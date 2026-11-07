# One-million-key recovery: measured

Status: **PASS. Recovery of 1,000,000 keys takes 0.55–0.80 s, under the 2 s limit, in every layout measured.** Measured 2026-11-07 on the hardware below. All numbers are warm-cache. Cold-cache was **NOT TESTED** (see "Limits").

## Environment

| Item | Value |
|---|---|
| CPU | AMD Ryzen 7 5800H (8 cores, 16 logical CPUs) |
| RAM | 32 GB |
| Disk | WD PC SN810 NVMe SSD (internal) |
| OS | Windows 11 Home 10.0.26200 |
| Go | go1.27.3 windows/amd64 |
| Build | `go build -o . ./src/cmd/kvdb` (default flags, no profiling) |

## What is timed

`recovery_ms` is the engine's own timer around the full `kvdb.Open` path: read the superblock, scan and checksum-verify every frame (CRC32C at all levels), apply each entry to the in-memory index, and apply the tail rules. The CLI prints it from `kvdb open <file>`. "Wall" is the whole fresh process, start to exit, including process start-up and lock-file handling.

Every run is a **fresh process** opening the file from disk; nothing is shared between runs except the OS file cache.

## Results

### 1. Worst case: one frame per key (1,000,000 frames)

Each key was its own committed PUT frame. That is the layout a client produces with 1M separate `put` calls, and it is the slowest to recover, because recovery checks one frame header and one payload checksum per key.

The file was written with the engine's own frame encoder (`format.WriteFrame`, the code path PUT uses) by a throwaway generator, deleted afterwards. That avoided 1M fsyncs; the bytes are the same as 1M real PUTs. `kvdb check` then verified it: `1000000 keys, 1000000 frames, 64888922 bytes … OK`. Spot reads returned `key0000000 → value-0` and `key0999999 → value-999999`.

| Run | recovery_ms | wall ms |
|---|---|---|
| 1 | 800.5 | 873 |
| 2 | 766.2 | 837 |
| 3 | 781.0 | 858 |
| 4 | 777.7 | 858 |
| 5 | 775.6 | 848 |

File: 64,888,922 bytes (65 MB). Keys `key0000000`–`key0999999` (10 bytes), values `value-<n>` (7–12 bytes).

### 2. Batched: 100 frames of 10,000 keys (CLI)

Loaded with 100 real `kvdb batch` invocations of 10,000 ops each (28 s in total, each batch fsynced before its ACK). File: 53,392,122 bytes, 100 frames. `kvdb check`: OK, 1,000,000 keys.

Five fresh-process `kvdb open` runs: recovery_ms **565–578**, wall **634–645 ms**.

### 3. In-process benchmark (`kvdb-perf --only recovery`)

Real `kvdb.Open` timed in-process, 3 runs each; data loaded in 10,000-op batches.

| Keys | File size | Median open | Min open |
|---|---|---|---|
| 100,000 | 12.3 MB | 35.4 ms | 33.3 ms |
| 1,000,000 | 123 MB | 557.9 ms | 547.1 ms |
| 2,000,000 | 246 MB | 1,206 ms | 1,199 ms |

Recovery time grows linearly. Even 2M keys (twice the target, with larger values) stay under 2 s.

## How a judge can reproduce it

```bash
go build -o . ./src/cmd/kvdb ./src/cmd/kvdb-perf
./kvdb-perf --only recovery          # in-process: 100k / 1M / 2M keys
```

To try the CLI path, write 100 batch files of 10,000 ops each (`kvdb batch <db> <file>` takes a file, one `put k v` per line), apply them, then time `open`:

```bash
for b in $(seq 0 99); do
  seq 0 9999 | sed "s/.*/put k$b-& v&/" > b.txt
  ./kvdb batch m1.kv b.txt >/dev/null
done
./kvdb open m1.kv          # prints keys=1000000 recovery_ms=... size=...
```

This exact loop was rehearsed: 37 s to load, then `keys=1000000 recovery_ms=548.120 size=23681232`. `kvdb-perf` is the quicker path.

## Limits (stated honestly)

* **Cold cache: NOT TESTED.** Flushing the Windows file cache needs administrator tools (RAMMap or a reboot), which were not used. In every run above, the file had just been written or read, so the OS cache held it. For a rough cold-cache estimate: reading 65 MB from this NVMe drive takes well under 0.1 s, so a cold open should stay below 2 s, but that is an estimate, not a measurement. On a slow spinning disk or a network drive, the result could differ.
* **One machine.** The judge's laptop may be slower. There is about 2.5× headroom in the worst case (0.80 s against 2 s).
* **Memory.** The in-memory index maps each live key to `{offset, len}` in the file; values stay on disk and are read on GET (DESIGN.md, index row of the structures table). 1M small keys fit comfortably.
