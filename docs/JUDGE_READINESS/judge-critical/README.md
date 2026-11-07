# Judge-critical test cases (8)

All 8 cases reuse the official scenario suite (`src/scenarios`); no duplicate tests were written. `run.sh` runs exactly these 16 scenarios on disposable temp databases. It records the real output in `runs/<timestamp>/`, never overwriting an earlier run:
* `run.log`: full `go test -v` output;
* `results.md`, `results.json`: per-scenario method, measurements and duration;
* `meta.json`: command, exit code, git HEAD.

It then copies the JSON to `frontend/src/data/judge-critical-results.json` for the dashboard's **Judge Readiness** page (`/judge-readiness`).

```bash
bash docs/JUDGE_READINESS/judge-critical/run.sh                     # about 1 minute
KVDB_SCENARIOS=full bash docs/JUDGE_READINESS/judge-critical/run.sh # full iteration counts
```

| # | Case | Scenarios | Kind of test |
|---|---|---|---|
| 1 | Torn writes | A2 | simulated crash (faultfs) |
| 2 | Batch atomicity | B2, B6 | simulated crash; B6 also real kills (torture large-batch) |
| 3 | Compaction crashes | C3, C4 | simulated crash (real-kill evidence: COMPACTION_SHRINK_TEST.md) |
| 4 | Interrupted recovery | C1, C2, F2 | simulated crash; F2 also real kills during `kvdb open` |
| 5 | Crash after fsync, before ACK | A4 | simulated crash (CrashAfter on Sync) |
| 6 | Concurrent writers | D2 | real process kills (torture same-key) |
| 7 | 1M-key recovery < 2 s | F3 | real process kill, timed `kvdb open` |
| 8 | Disk full | E1–E5 | real files with the max_bytes cap, injected ERROR_DISK_FULL, simulated crash, real kills (E3) |

**Simulated crash** means faultfs stops the real engine at one exact file operation, in-process, and drops its handles without cleanup. **Real process kill** means `TerminateProcess` on a real `kvdb` child. Neither is a power cut. A physically full disk and a physical power cut are **NOT TESTED**.

Latest run (2026-11-07 10:55, quick sizes): **16/16 scenarios PASS; all 8 cases PASS.** F3 recovered 1,000,000 acknowledged keys in 531 ms (555 ms process wall time).
