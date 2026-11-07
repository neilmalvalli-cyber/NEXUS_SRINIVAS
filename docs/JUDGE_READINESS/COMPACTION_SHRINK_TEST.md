# Compaction shrinks the file: measured

Status: **PASS.** A 103 MB database with 90% of its keys deleted shrank to 10.3 MB, and every remaining key and value was intact. Process kills during compaction lost nothing. Measured 2026-11-07 with the real `kvdb` CLI, on disposable files in the session scratch folder. Hardware is as in ONE_MILLION_KEY_RECOVERY.md.

## Test 1: delete 90%, then compact

**Setup.** 100,000 keys (`key000000`–`key099999`), each with a value of about 1 KB (`v<n>-` followed by 1,000 `x`), loaded as 10 `kvdb batch` files of 10,000 puts.

| Step | File size | `kvdb check` |
|---|---|---|
| After load | 102,800,352 B (103 MB) | 100,000 keys, 10 frames, OK |
| Delete batches 0–4 (45,000 deletes) | 102,980,384 → 103,700,512 B | (file grows: deletes are appended records) |
| Delete batch 5 | **47,288,416 B** | automatic compaction ran: the file was ≥ 4 MiB and more than 2× its compacted size |
| Delete batches 6–7 | 47,468,448 → 47,648,480 B | |
| Delete batch 8 | **19,532,192 B** | automatic compaction again |
| Delete batch 9 (90,000 deletes in total) | 19,712,224 B | 10,000 keys, 6 frames, OK |
| Explicit compaction (`w1 c1 compact` via `kvdb serve`, replied `ACK c1`) | **10,280,128 B (10.3 MB)** | 10,000 keys, 3 frames, 10,280,000 live bytes, OK |

The final file is 10.0% of its peak size, and only 128 bytes larger than the live data (superblock plus frame headers).

**Verification after compaction**
* `kvdb dump` printed exactly 10,000 keys.
* `key000000` and `key099990` (kept) returned their full values.
* `key000001` and `key099999` (deleted) returned `NOTFOUND`.

## Test 2: kill the process during compaction

**Setup.** 100,000 × 1 KB keys, then 40% deleted (keys whose number mod 5 is 0 or 1), as 20 frames. That leaves 103,600,672 B on disk for 61,680,000 live bytes, so automatic compaction does not trigger (the file is under 2× its compacted size). For each run, a fresh copy was made, `kvdb serve` received `compact`, and the process was killed with `taskkill /F` after a delay. The file was then reopened.

| Kill after | `ACK` seen | Leftover before reopen | Size after reopen | check | dump |
|---|---|---|---|---|---|
| 20 ms | no | `t.kv.compact` (unfinished) | 103,600,672 (old file kept) | OK | 60,000 |
| 60 ms | no | `t.kv.compact` | 103,600,672 (old file kept) | OK | 60,000 |
| 120 ms | no | `t.kv.compact` | 103,600,672 (old file kept) | OK | 60,000 |
| 250 ms | no | `t.kv.old` (swap had happened) | 61,680,512 (new file kept) | OK | 60,000 |
| 500 ms | yes | none | 61,680,512 | OK | 60,000 |
| 1000 ms | yes | none | 61,680,512 | OK | 60,000 |

**Repeat check** of the 60 ms and 250 ms points, with deleted-key checks:
* Open deleted the leftover `t.kv.compact`, leaving only `t.kv` and the `.lock` file.
* `key000000`, `key000001` and `key000005` (deleted) were `NOTFOUND`; `key099999` (live) was intact.
* A scan of all 60,000 dumped keys found 0 deleted keys.

In every case the database held exactly the 60,000 live keys and none of the deleted ones. It was either the old file (compaction had not committed) or the new compacted file (compaction had committed), never a mix. That is the documented A1–A7 authority rule (DESIGN §11).

Systematic coverage of compaction crashes: `./torture --mode compaction` (real kills at random points; see CRASH_SCENARIO_RESULTS.md) and the storage tests that crash at each compaction step.

## Notes

* Deletes make the file **grow** until compaction runs, because the log is append-only. That is by design.
* Compaction never makes the file larger; when nothing can be reclaimed it does nothing (`kvdb.DB.Compact` doc comment).
* A kill is a process crash, not a power cut. Power loss during compaction is covered only by the simulated Power-Cut model (POWER_FAILURE_MODEL.md).

## Reproduce

```bash
go build -o . ./src/cmd/kvdb
PAD=$(printf 'x%.0s' $(seq 1 1000))
for b in $(seq 0 9); do
  awk -v b=$b -v p=$PAD 'BEGIN{for(i=0;i<10000;i++){k=b*10000+i; printf "put key%06d v%06d-%s\n",k,k,p}}' > b.txt
  ./kvdb batch s.kv b.txt
done
ls -l s.kv                                     # ~103 MB
for b in $(seq 0 9); do
  awk -v b=$b 'BEGIN{for(i=0;i<10000;i++){k=b*10000+i; if(k%10) printf "del key%06d\n",k}}' > d.txt
  ./kvdb batch s.kv d.txt
done
printf 'w1 c1 compact\n' | ./kvdb serve s.kv   # ACK c1
ls -l s.kv                                     # ~10.3 MB
./kvdb check s.kv
```

One batch holds at most 10,000 operations. A larger batch is refused as a whole (`ERROR (too-large)`) and nothing is applied, so keep each delete file under 10,000 lines.
