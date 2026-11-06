# Validation run (Prompt 10), measured 2026-11-06

Every file here was written by one run of
[run-validation.sh](run-validation.sh) against the current working tree
(Prompt 10 changes included). It started at 19:11 IST and ended at 19:58 IST
with `script exit 0`.

Machine: Windows 11 Home 10.0.26200, AMD Ryzen 7 5800H (16 threads), 32 GB
RAM, NVMe SSD, Defender real-time protection on, Go 1.27.3 windows/amd64.

To reproduce, run this from the repository root in Git Bash. It takes about
50 minutes and overwrites the logs here.

```bash
bash docs/results/validation/run-validation.sh
```

| File | What | Result |
|---|---|---|
| [00-console.log](00-console.log) | summary of every stage | `script exit 0` |
| [01-static-and-unit.log](01-static-and-unit.log) | gofmt, vet ×4 OSes, build, `go test -count=1 ./...` | clean; all ok |
| [02-crosscheck.log](02-crosscheck.log) | `crosscheck/run.sh` against the independent implementation | 9/9 PASS |
| [03-targeted-tests.log](03-targeted-tests.log) | Windows move tests, `recovery.Inspect`, `check --no-repair`, deterministic compaction | ok |
| [04-scenarios-full.log](04-scenarios-full.log), [SCENARIO_RESULTS.md](SCENARIO_RESULTS.md) | 32 official scenarios, `KVDB_SCENARIOS=full` | 32/32 PASS, 909 s |
| [05-perf-1M-recovery.log](05-perf-1M-recovery.log) | open of 1,000,000 keys | 467 ms |
| [06-acceptance-1M.log](06-acceptance-1M.log), [acceptance-1M-put-delete.txt](acceptance-1M-put-delete.txt) | 1M synced PUTs, then open, 1M DELETEs, then compaction, then 10k writes and a reopen | PASS: open 661 ms; 101.7 MB → 32 bytes |
| `torture-*.log` | 8 torture runs (seeds in the file names) | all PASS: 500/500 and 7 × 100/100 |

JSON reports and oracle logs of the torture runs are under
`torture-runs/validation-*` (ignored by git).

**Not covered by this run:**

* `go test -race` (no C compiler on this machine);
* power-cut tests.

Torture Engine 2.0 (`nexus-torture`) was validated later, separately:
[../../TORTURE_RESULTS.md](../../TORTURE_RESULTS.md).

In every real-kill torture run, recovery never had to cut a torn tail
(`torn-tails=0`). Torn frames are covered by the fault-injection scenarios.
