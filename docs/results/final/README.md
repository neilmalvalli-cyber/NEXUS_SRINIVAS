# Final validation (Prompt 12/13), 2026-11-06 22:15–23:13 IST

Machine: Windows 11 Home Single Language build 26200, AMD Ryzen 7 5800H
(16 logical CPUs), 32 GB RAM, NVMe SSD, Windows Defender real-time protection
on, Go 1.27.3 windows/amd64. Code: commit `8058c2d` **plus the uncommitted
working tree** (listed in [01-environment.log](01-environment.log)). Engine
source (`src/kvdb`, `src/internal`, `src/cmd/kvdb`) last changed 19:06:59;
nothing in it changed during or after this run.

How it ran:

1. [run-final.sh](run-final.sh) ran steps 01–08 and the Torture Engine 2.0
   default run. It was then **stopped on purpose** at 22:37:15, before its
   remaining steps. Those steps repeat evidence already recorded for the same
   engine code: the 11 other torture modes (earlier tester build, 21:30), the
   full-size scenario run and the 1,000,000 PUT/DELETE acceptance
   ([../validation/](../validation/README.md), 19:11–19:58). The batch10 run
   it had just started was stopped before its first iteration finished;
   [torture/batch10-100-seed2026.log](torture/batch10-100-seed2026.log) holds
   only its two header lines and is not a result.
2. [run-targeted.sh](run-targeted.sh) then ran mutation testing, the
   detection rate, reproducibility, all 32 scenarios (quick) and the
   benchmark, stopping at the first failure (none occurred).

Every step and exit code: [00-steps.log](00-steps.log).

## Results

| # | Step | Command (abridged) | Exit | Result | Log |
|---|---|---|---|---|---|
| 01 | Environment | `go version; git status` | — | recorded | [01](01-environment.log) |
| 02 | Clean build | `go build -a` of kvdb, kvdb-bench, nexus-torture, kvdb-mutant into an empty directory | 0 | ok | [02](02-clean-build.log) |
| 03 | Static checks | `gofmt -l`; `go vet` for windows, linux, darwin, freebsd; `go build ./...` incl. linux/arm64 | 0 | gofmt clean, vet ok ×4, build ok | [03](03-static.log) |
| 04 | Unit and integration tests | `go test -count=1 ./...` | 0 | 18 packages ok, 0 failed | [04](04-go-test.log) |
| 05 | Cross-check (independent implementation) | `crosscheck` | 0 | **9 checks, 0 failed** | [05](05-crosscheck.log) |
| 06 | Targeted: compaction, disk full / size cap, read-only check, Windows moves | `go test -v -run 'Compact\|DiskFull\|MaxBytes\|Full\|Inspect\|CheckNoRepair\|Move\|KillDuringCompacting'` | 0 | 375 subtests PASS, 0 FAIL | [06](06-targeted.log) |
| 07 | 1,000,000-key recovery | `KVDB_PERF=1 go test ./src/internal/recovery -run TestOpenMillionKeysTiming` | 0 | **open of 1,000,000 keys (37.0 MB): 474 ms** (limit 2 s) | [07](07-recovery-1M.log) |
| 08 | 32 official scenarios, Go fault-injection suite, full mode | `KVDB_SCENARIOS=full go test ./src/scenarios -run TestGroup` | 0 | **32 / 32 PASS** (918 s; A 91 s, B 82 s, C 84 s, D 256 s, E 67 s, F 336 s) | [08](08-scenarios-gotest-full.log), [table](SCENARIO_RESULTS_gotest.md) |
| T1 | Torture 2.0, judge invocation | `nexus-torture run --mode default --seed 12345 --iterations 500` | 0 | **PASS 500/500**, 0 inconclusive; 148,100 acked writes, all verified after restart; 0 batch-atomicity, 0 integrity, 0 recovery failures | [log](torture/default-500-seed12345.log), [summary](torture/default-500-seed12345.summary.json) |
| T2 | Mutation testing | `nexus-torture mutants --iterations 40 --seed 777` | 0 | **all 6 mutations detected at iteration 1; control passed** | [log](torture/mutants.log) |
| T3 | Detection rate | 20 iterations per mutation, `--keep-going`, seed 4040 | 0 | 20/20 ×4; partial-batch 13/20; bad-recovery 5 of 7 valid iterations (13 invalid, environment) | [log](torture/mutation-detection-rate.log) |
| T4 | Reproducibility | seed 31337 twice + replay of a recorded failure | 0 | both PASS 20/20; 14,883 requests compared, **0 mismatches**; replay reproduced seed, kill time and violation kind | [log](torture/reproducibility.log) |
| T5 | 32 scenarios against the real executable | `nexus-torture scenarios` (quick sizes) | 0 | **32 / 32 PASS**, 0 inconclusive, 1,385 kill points / crash states, 267 s | [log](torture/scenarios-quick.log), [json](torture/scenarios-quick.json) |
| 09 | Benchmark | `kvdb-bench --n 100000 --value-size 100 --batch-size 1000 --runs 3 --seed 1` | 0 | see below | [log](09-bench.log), [BENCHMARK.md](BENCHMARK.md), [bench.json](bench.json) |

Explanations and the detection-rate details: [../../TORTURE_RESULTS.md](../../TORTURE_RESULTS.md).

## Benchmark (median of 3 runs)

Every put, delete and batch is synced to disk before it returns (one
`FlushFileBuffers` per call). In-process API, 100,000 keys, 100-byte values,
batches of 1,000. One machine; numbers depend on the disk. No comparison
with other databases is made or implied.

| Phase | Ops | Seconds | Ops/s | p50 µs | p99 µs | Auto compactions | File before → after (bytes) |
|---|---|---|---|---|---|---|---|
| put | 100,000 | 37.05 | 2,699 | 520 | 1,064 | 0 | → 15,500,032 |
| reopen (recovery of 100,000 keys) | — | 0.045 | — | — | — | 0 | 15,500,032 |
| get | 100,000 | 0.60 | 165,652 | — | — | 0 | — |
| batch (100 × 1,000 ops) | 100,000 | 0.55 | 180,946 | 1,034 per batch | 460,836 per batch | 1 | → 15,498,960 |
| compact (100,000 live keys) | — | 0.46 | — | — | — | — | 15,498,960 → 12,300,128 |
| delete | 100,000 | 37.49 | 2,668 | 520 | 1,266 | 3 | → 3,680,926 |
| compact (empty) | — | 0.006 | — | — | — | — | 3,680,926 → 32 |

The batch p99 is the one batch that triggered an automatic compaction. Get
latency is not reported per call because it is below the timer resolution.

## What is not covered by this run (and where it is)

| Not run here | Covered by |
|---|---|
| 11 non-default torture modes with the final tester build | earlier tester build, same engine, all PASS: [torture/earlier-tester-build-2130/](torture/earlier-tester-build-2130/) |
| `nexus-torture scenarios --full` with the final build | Go suite in full mode (step 08); earlier build passed A1–C1 at full size |
| 1,000,000 PUT + 1,000,000 DELETE acceptance | [../validation/](../validation/README.md), same engine code |
| Race detector | not available (no cgo on this machine) |
| Power loss, Linux/macOS execution | not tested |
