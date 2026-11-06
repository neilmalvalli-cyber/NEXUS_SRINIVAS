# Performance optimization (Prompt 19)

What was measured, what was changed, what was rejected, and what it cost.
Durability, crash consistency and the on-disk format were fixed constraints:
every acknowledged write is still fsynced before it returns, and the file
format is byte-for-byte unchanged (no format version bump, no migration).

All numbers below are **recorded** on one machine (AMD Ryzen 7 5800H, 16
logical CPUs, Windows 11, internal disk of unrecorded type, Go 1.27.3) with
the same `kvdb-perf` workload for every step. They are machine-dependent and
are not a comparison with any other database. The raw files are in
[results/optimization/](results/optimization/), and the dashboard's Benchmark
page renders [history.json](results/optimization/history.json), which is
generated from them.

## 1. Method

* **Tool.** `src/cmd/kvdb-perf` drives the public `kvdb` API in-process. It
  measures throughput and latency percentiles for 1/4/16 concurrent writers,
  batch and delete, GET with 1 and 8 threads, a mixed load (4 readers + 4
  writers), heap bytes per key at 1M keys, open (recovery) time at 0.1M/1M/2M
  keys, and compaction of a 50%-dead 100k-key file with two readers running
  (compaction time plus the longest reader wait). `--profiles DIR` writes
  CPU, heap, mutex and block profiles per phase.
* **Baseline.** The engine as of commit `7a1f536`, built as a separate
  binary, with profiles.
* **One change at a time.** After each change: `go test ./...`, then the same
  `kvdb-perf` run, then a comparison. A change that did not help was not kept.
* **Validation of the final engine.** `go test ./...`, the torture runs listed
  in §6, and the Power-Cut Explorer ([POWER_CUT_EXPLORER.md](POWER_CUT_EXPLORER.md)).

### What the baseline profiles showed

| Profile | Finding |
|---|---|
| `writers-16` CPU | 96% of samples in `fileio.osFile.Sync` (the syscall). A writer's cost is the fsync, and 16 writers each paid their own fsync one after another. |
| `mixed` mutex | 99% of the contention was writers releasing the handle's `RWMutex` held across write + fsync + index update. Readers queued behind every fsync. |
| `get-8` block | 79% of blocked time in `internal/poll.(*fdMutex).rwlock` under `ReadAt`: on Windows, positioned reads on one `*os.File` serialize. |
| `recovery-1000000` CPU | Dominated by Go map hashing and insert (index rebuild) and CRC32C; the scan already reads sequentially through a 4 MiB buffer. |
| `compaction` block | Readers were blocked for the whole copy phase (≈ 0.5 s). |

## 2. The six areas evaluated

| Area | Decision | Why |
|---|---|---|
| **A. Group commit** | **Done** | The fsync is the cost. Commits that arrive while another is syncing now share the next frame and its single fsync. |
| **B. Locking** | **Done** | Split the handle lock so readers do not wait for fsync or for compaction's copy. |
| **C. Index memory / read path** | Read path **done** (read-handle pool); index packing **rejected** | Parallel GETs serialized on one Windows file handle. Packing offset+size into one word saves ~8 B/key but caps file size and fixes no measured bottleneck. |
| **D. Serialization** | **Rejected** | Any change to how frames are encoded is a format change. The encoder already stages a frame and writes it with one `WriteAt`. |
| **E. Recovery** | **No change** | One sequential buffered pass with 2 allocations per key (the key string and the map entry). The remaining cost is the hash map itself. |
| **F. Compaction** | Improved **through B**; no algorithm change | Readers keep running during the copy. Compaction itself takes longer under read load (trade-off, §4). |

### A. Group commit (`storage/log.go`: `Commit`, `runGroup`)

* Each `Commit` enqueues its request (under a small queue mutex), then
  competes for the writer lock `wmu`. Whoever gets `wmu` takes **every queued
  request**, merges consecutive requests into one frame (bounded by
  `format.MaxFrameOps` operations and `GroupPayloadCap` = 8 MiB of payload),
  writes it, fsyncs once, publishes the index changes and marks each request
  done. Followers whose request was already committed return its result.
* **No timer.** A lone writer commits immediately; grouping happens only when
  commits pile up behind an fsync that is already running.
* **Durability is unchanged.** A caller returns only after the fsync that
  covers its operations. Operations merged into one frame are durable together
  or not at all: recovery applies frames whole, so a merged frame is exactly as
  atomic as a batch.
* **Admission.** A merged commit must be a plain append. If it would need a
  compacting commit or would exceed `max_bytes`, the group is split
  (`errSplit`) and each request is admitted on its own, so `ErrFull` is still
  decided per request (test: `TestGroupCommitSplitsUnderMaxBytes`).
* **Format.** A merged frame is an ordinary frame with `op_count > 1`, which
  the format and recovery already accepted (batches). No new field.

### B. Lock split (`wmu` / `mu`)

* `wmu` (mutex) serializes everything that changes the database: commits,
  compaction and close. `mu` (RWMutex) protects what readers see. Lock order:
  `wmu` before `mu`.
* A commit holds `wmu` through write and fsync and takes `mu` exclusively only
  for the in-memory publish (index update, counters). Compaction holds `wmu`
  for the whole copy and takes `mu` only for the final swap.
* Invariant I1 (at most one unsynced frame, always the last) is kept: only the
  `wmu` holder writes.

### C. Read-handle pool (`storage/readpool.go`)

* Up to 4 extra read-only handles on the data file, handed out to concurrent
  GETs from a channel. They are closed and reopened around compaction's swap,
  which holds both `wmu` and `mu`, so no reader holds a handle to a replaced
  file.

## 3. Before / after (recorded)

Baseline = commit `7a1f536`; final = A + B + C. The intermediate steps are in
`history.json` and on the Benchmark page. GET and compaction baselines come
from the second baseline run (`02-baseline-get-compaction.json`), because the
first run's compaction workload was flawed and its GET numbers varied widely
(64,255/s and 138,108/s on the first run).

| Metric | Baseline | B | B+C | Final (A+B+C) |
|---|---:|---:|---:|---:|
| PUT, 1 writer (ops/s) | 2,585 | 2,646 | — | 2,636 |
| PUT, 4 writers (ops/s) | 2,606 | 2,603 | — | **4,659** |
| PUT, 4 writers p50 (µs) | 1,570 | 1,569 | — | **556** |
| PUT, 16 writers (ops/s) | 2,442 | 2,613 | — | **12,426** |
| PUT, 16 writers p50 / p99 (µs) | 6,358 / 8,998 | 6,009 / 7,546 | — | **1,203 / 3,149** |
| BATCH of 100 (ops/s) | 201,271 | — | — | 198,121 |
| DELETE, 1 writer (ops/s) | 2,565 | — | — | 2,664 |
| GET, 1 thread (ops/s) | 157,629 | — | 158,617 | 150,680 |
| GET, 8 threads (ops/s) | 108,661 | — | 316,043 | **312,699** |
| Allocations per GET | 2.00 | — | 2.00 | 2.01 |
| Mixed: GET (ops/s) | 7,842 | 10,556 | 166,781 | **231,568** |
| Mixed: GET p99 / max (µs) | 1,561 / 20,633 | 1,637 / 4,506 | 1,068 / 1,972 | **1,002 / 1,960** |
| Mixed: PUT (ops/s) | 1,953 | 2,167 | 2,023 | **4,366** |
| Compaction, 2 readers running (ms) | 502 | 3,080 | 1,057 | 848 (slower, §4) |
| Longest reader wait during compaction (µs) | 501,739 | 36,032 | 34,980 | **37,223** |
| Index heap per key, 1M keys (B) | 99.85 | — | — | 99.81 |
| Open (recovery), 1M keys (ms) | 580 | — | — | 512 |
| Open (recovery), 2M keys (ms) | 1,242 | — | — | 1,107 |

How to read it:

* Single-writer PUT and DELETE are unchanged (within variance). They are bound
  by one fsync each, and nothing here may remove that fsync.
* The recovery code was not changed, so its 10% difference is run-to-run
  variance, not an improvement. The same goes for single-thread GET (±5%).
  The dashboard greys out any change within ±5%.
* B alone made compaction much slower (3,080 ms): readers that were no longer
  blocked all competed for the single file handle. C (the handle pool) brought
  most of that back.

## 4. Trade-offs

* **Compaction under concurrent reads takes longer** (502 → 848 ms), because
  readers now share the disk and CPU during the copy instead of waiting. In
  exchange, the longest reader wait fell from ~502 ms to ~37 ms. Compaction
  without concurrent readers was not measured separately.
* **Group atomicity.** Commits merged into one frame survive a crash together
  or not at all. Since no merged caller has been acknowledged before the fsync,
  this never loses an acknowledged write. It does mean that two independent
  unacknowledged writes can disappear together.
* **Four more open handles** per database (read-only).

## 5. Rejected ideas

| Idea | Reason |
|---|---|
| Packed index location (offset+size in one `uint64`) | Saves ~8 B/key but caps the file at ~8 TiB and changes no measured bottleneck. |
| Faster recovery (parallel scan, hint files) | Hint files or checkpoints are a format change; a parallel scan would complicate the strictly sequential rules C0–C8. Considered, not implemented or measured. |
| Serialization changes | Would change the on-disk format. |
| Timer-based group commit / delayed fsync | Adds latency for a lone writer, or weakens durability. Not allowed. |
| Skipping the per-GET entry CRC | Would weaken corruption detection on reads. Considered, not attempted. |

## 6. Validation of the optimized engine

* `go test ./...` passed after every change. New tests:
  `storage/concurrency_test.go` (6 writers + 4 readers with batch-atomicity and
  reopen-equality checks; group commit under `max_bytes`).
* Torture with the optimized binaries, logs in
  [results/optimization/](results/optimization/):
  * `torture-default-300-seed12345.log`: 300 iterations, 144,671 acknowledged
    writes verified, PASS;
  * `torture-compaction-100-seed7.log`, `torture-large-batch-60-seed13.log`,
    `torture-disk-full-60-seed11.log`: PASS;
  * `torture-mutants.log`: every planted bug detected, control passed.
* Power-Cut Explorer default run (`powercut-default-seed1.json`): 1,278 modeled
  power-loss images from 291 crash points recovered correctly, and 6/6
  negative controls were detected. That is a finite sample, not a proof.
* The race detector was **not** run: it needs cgo (gcc), which is not
  installed on the measurement machine (`CGO_ENABLED=0`). Concurrency is
  covered by the stress tests and torture runs above instead.

## 7. Reproduce

```
go build -o kvdb-perf ./src/cmd/kvdb-perf
kvdb-perf --json perf.json [--profiles prof/] [--only writers,get,mixed] [--quick]
```

From the dashboard (Benchmark → *Run kvdb-perf now*), runs use `--quick`
sizes, are labelled LIVE, and are never merged with the recorded history.
