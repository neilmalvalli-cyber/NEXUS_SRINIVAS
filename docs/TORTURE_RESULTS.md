# NEXUS Torture Engine 2.0 — measured results

Everything below was produced by commands that actually ran on 2026-11-06,
on one machine: Windows 11 Home 10.0.26200, AMD Ryzen 7 5800H (8 cores / 16
threads), 32 GB RAM, NVMe SSD, Windows Defender real-time protection **on**,
Go 1.27.3. The logs and JSON summaries are in
[results/final/torture/](results/final/torture/). Nothing is an estimate.

Two kinds of evidence are kept apart:

* **Current tester build**: `nexus-torture` built at 22:15 from the final
  code (`go build -a`, in `torture-runs/final/bin`). Runs from 22:34 to
  23:13 by [run-final.sh](results/final/run-final.sh) and
  [run-targeted.sh](results/final/run-targeted.sh).
* **Earlier tester build (21:30)**: the same `kvdb` engine (engine source
  unchanged since 19:06:59), but a `nexus-torture` from before the last
  tester fixes (verification counters, the `--keep-going` baseline fix, the
  rewritten C6 and A7 scenarios). These runs are kept as evidence about the
  engine. Their summaries were copied unchanged to
  [results/final/torture/earlier-tester-build-2130/](results/final/torture/earlier-tester-build-2130/).

A run is PASS only when the oracle verified every iteration. INCONCLUSIVE is
counted separately and is never a pass. No run below had an inconclusive
iteration.

## 1. Judge invocation — current tester build

`nexus-torture run --mode default --seed 12345 --iterations 500`
([log](results/final/torture/default-500-seed12345.log),
[summary.json](results/final/torture/default-500-seed12345.summary.json)):

```
RESULT: PASS  mode=default seed=12345  passed=500 failed=0 inconclusive=0 of 500  acked=157731 rejected=0 unknown=1351 gets=39609  kills-mid-write=488 compact-in-flight=321 torn-tails=0 interrupted-compactions=7 recovery-kills=0/0 closes=0 kills-in-close=0
VERIFY: attempted=198834 acknowledged=197340 (writes 148100) cleanly-rejected=0 unknown=1351  acked-writes-verified-after-restart=148100 reads-verified=39609  batch-atomicity-violations=0 data-integrity-violations=0 recovery-failures=0 protocol/liveness-failures=0 oracle-inconclusive=0
PERF: 180.5s total, 2.77 iterations/s, restart 11.6 ms avg / 15.8 max, recovery 1.44 ms avg / 3.22 max, oracle 0.76 ms avg / 3.6 max, verify 35.5 ms avg, serve peak 24.0 MiB, tester heap peak 5.5 MiB
```

| Counter | Value |
|---|---|
| Operations attempted | 198,834 |
| Acknowledged (all kinds) | 197,340 |
| Acknowledged data writes (put / delete / batch) | 148,100 |
| Acknowledged writes verified present after restart | **148,100 (100 %)** |
| Reads verified against the oracle | 39,609 |
| Cleanly rejected | 0 |
| Unknown outcome (in flight at the kill; accepted only as all-or-nothing) | 1,351 |
| Batch atomicity violations | **0** |
| Data integrity violations | **0** |
| Recovery failures | **0** |
| Oracle inconclusive | **0** |
| Kills with a write in flight | 488 of 500 |
| Kills with a compaction in flight | 321 |
| Recoveries that completed an interrupted compaction | 7 |

The RESULT line's `acked=157,731` counts acknowledged writes **and**
compactions; the VERIFY line separates data writes (148,100) from reads, so
`acknowledged = 148,100 writes + 39,609 reads + 9,631 compactions`.

## 2. The 32 official scenarios against the real executable — current build

`nexus-torture scenarios` (quick sizes)
([log](results/final/torture/scenarios-quick.log),
[scenarios-quick.json](results/final/torture/scenarios-quick.json),
[table](results/final/torture/SCENARIOS-quick.md)):

```
SCENARIOS RESULT: PASS  32 passed, 0 failed, 0 inconclusive/unsupported, of 32  (267s)
```

1,385 kill points or crash states in total, every one checked. A7 (1 MiB
put) and C6 (kill during close) are the rewritten versions. Quick sizes use
smaller databases than the official sizes (for example F3 and B6 do not use
1,000,000 keys here).

**Full sizes (`scenarios --full`) were not re-run with the current build.**
The 1,000,000-key and 500-kill sizes are covered for the same engine by the
Go suite in full mode (section 6) and by the earlier tester build, which
passed A1–C1 at full size before that run was stopped (section 5).

## 3. Mutation testing — current build

Six deliberately broken versions of the database are built as a separate
binary, `kvdb-mutant`. The official `kvdb` binary is never modified.

First detection, `nexus-torture mutants --iterations 40 --seed 777`
([log](results/final/torture/mutants.log),
[mutation-report.json](results/final/torture/mutation-report.json)):

```
MUTATION RESULT: every mutation detected, control passed
  none             as expected  iteration=0   kind=
  ack-before-sync  as expected  iteration=1   kind=acknowledged-write-lost
  drop-acked       as expected  iteration=1   kind=invalid-read
  partial-batch    as expected  iteration=1   kind=partial-batch
  wrong-unknown    as expected  iteration=1   kind=rejected-write-visible
  bad-recovery     as expected  iteration=1   kind=acknowledged-write-lost
  corrupt-value    as expected  iteration=1   kind=value-never-written
```

Detection rate: 20 iterations per mutation, `--keep-going`, seed 4040
([log](results/final/torture/mutation-detection-rate.log)):

| Mutation | Iterations failed (= detected) | Notes |
|---|---|---|
| ack-before-sync (ACK before fsync) | **20 / 20** | |
| drop-acked (an acknowledged write is lost) | **20 / 20** | |
| wrong-unknown (a rejected write is applied) | **20 / 20** | |
| corrupt-value (every 6th value has one byte changed) | **20 / 20** | |
| partial-batch (batch written as separate frames) | **13 / 20** | the fault is only visible if the kill lands *between* the frames of an in-flight batch; the 7 passing iterations had a batch in flight but the kill fell before or after all of its frames, which leaves a state a correct database could also produce. An earlier run with the same command measured 19 / 20; kill timing is scheduler-dependent |
| bad-recovery (recovery drops the last acknowledged put) | **5 of 7 valid iterations** | iterations 4–16 are **invalid**: see below |

**Invalid `bad-recovery` iterations 4–16 (environment, mutant-only).** From
iteration 4 every "open after crash" of the mutant timed out after 2 minutes,
and the next serve exited after 3 requests. Investigation (22:55–23:05):
opening the mutant's side file `db.kv.mutant-last` blocked for **every**
process on the machine (the mutant, Git's `ls`, and .NET `File.Open`), while
the same database bytes, copied elsewhere, opened in 60 ms with both the real
`kvdb` and the mutant. No `kvdb` or `kvdb-mutant` process from the earlier
iterations was alive. The file is written only by the mutant wrapper; the
real engine never creates or reads it. The stall cleared by itself around
23:00, after which iterations 17–20 ran normally. The most likely cause is
a file-system filter (Defender real-time protection was on) holding a file
that a killed process had just written. That is not proven, because no
handle-inspection tool was available. These 13 iterations are reported as
invalid and not counted as detections.

## 4. Reproducibility — current build

([log](results/final/torture/reproducibility.log))

| Check | Result |
|---|---|
| `run --seed 31337 --iterations 20`, twice | both **PASS 20/20** |
| Iteration seeds and kill times | identical in both runs |
| Request streams (60 writer streams, 14,883 requests compared) | **0 mismatches** |
| Replay of a recorded failure (mutant `corrupt-value`, iteration 5) with `--iteration 5 --from-db failures/iter-5/pre/db.kv` | same iteration seed (2731518401891682087), same kill time (143 ms), same violation kind (`value-never-written`); the affected key differs (k0042 vs k0021) because the mutant's every-6th-put counter depends on how the three writers interleave |

The workload is reproducible exactly. The kill lands at the same requested
time, but what the process had finished by then depends on the OS scheduler.

## 5. All 12 modes — earlier tester build (21:30), same engine

`nexus-torture run --mode M --seed 2026 --iterations 100` (default: seed
12345, 500 iterations), all PASS, 0 failed, 0 inconclusive. Summaries:
[earlier-tester-build-2130/](results/final/torture/earlier-tester-build-2130/).

| Mode | Seed | Result | Acked writes | Rejected (`full`) | Unknown | Gets verified | Notable | Time |
|---|---|---|---|---|---|---|---|---|
| `default` | 12345 | **PASS 500/500** | 142,844 | 0 | 1,365 | 36,038 | 335 kills with compaction in flight; 25 interrupted compactions resolved | 188 s |
| `batch10` | 2026 | **PASS 100/100** | 66,347 | 0 | 271 | 16,810 | 1 interrupted compaction resolved | 39 s |
| `large-values` | 2026 | **PASS 100/100** | 8,530 | 0 | 184 | 1,510 | 1 interrupted compaction resolved; 1 torn tail discarded | 88 s |
| `large-batch` | 2026 | **PASS 100/100** | 103,022 | 0 | 283 | 17,188 | — | 186 s |
| `concurrent-disjoint` | 2026 | **PASS 100/100** | 68,915 | 0 | 177 | 12,239 | 1 interrupted compaction resolved | 39 s |
| `same-key` | 2026 | **PASS 100/100** | 46,043 | 0 | 185 | 5,114 | 2 interrupted compactions resolved | 38 s |
| `readers` | 2026 | **PASS 100/100** | 87,179 | 0 | 98 | 240,679 | — | 87 s |
| `compaction` | 2026 | **PASS 100/100** | 36,781 | 0 | 288 | 3,352 | 89 kills with compaction in flight; 6 interrupted compactions resolved | 84 s |
| `recovery-crash` | 2026 | **PASS 100/100** | 1,523 | 0 | 280 | 422 | 153 of 205 kills landed during recovery | 282 s |
| `disk-full` | 2026 | **PASS 100/100** | 11,554 | 5,558 | 299 | 916 | every rejected write verified absent | 63 s |
| `close-reopen` | 2026 | **PASS 100/100** | 28,348 | 0 | 137 | 7,097 | 47 graceful closes, 13 killed during close | 36 s |
| `mixed-long` | 2026 | **PASS 100/100** | 339,876 | 0 | 376 | 85,035 | 68 kills with compaction in flight | 529 s |

Performance of the tester in those runs:

| Mode | Iterations/s | Restart ms avg / max | Recovery ms avg / max | Oracle ms avg / max | serve peak MiB | tester heap peak MiB |
|---|---|---|---|---|---|---|
| `default` | 2.66 | 12.8 / 18.1 | 2.14 / 4.27 | 0.72 / 2.6 | 22.2 | 4.9 |
| `batch10` | 2.56 | 17.0 / 26.2 | 5.69 / 10.62 | 2.40 / 7.3 | 20.3 | 10.9 |
| `large-values` | 1.13 | 20.9 / 34.2 | 8.82 / 17.98 | 0.14 / 0.6 | 35.6 | 74.9 |
| `large-batch` | 0.54 | 30.2 / 43.0 | 18.41 / 29.72 | 206.99 / 583.6 | 29.4 | 255.1 |
| `concurrent-disjoint` | 2.54 | 17.8 / 28.1 | 6.52 / 13.76 | 1.10 / 2.8 | 17.1 | 9.6 |
| `same-key` | 2.62 | 15.3 / 20.9 | 3.28 / 5.89 | 1.76 / 5.9 | 19.4 | 8.7 |
| `readers` | 1.15 | 20.5 / 34.7 | 9.56 / 21.79 | 297.72 / 825.7 | 20.3 | 15.2 |
| `compaction` | 1.20 | 12.9 / 16.2 | 2.52 / 6.79 | 0.52 / 1.3 | 25.9 | 10.0 |
| `recovery-crash` | 0.35 | 69.1 / 76.7 | 55.89 / 63.85 | 134.72 / 158.7 | 60.1 | 68.0 |
| `disk-full` | 1.59 | 13.1 / 16.7 | 2.41 / 4.27 | 0.24 / 1.1 | 23.4 | 4.6 |
| `close-reopen` | 2.74 | 12.4 / 14.3 | 1.98 / 4.14 | 0.72 / 2.1 | 22.2 | 4.9 |
| `mixed-long` | 0.19 | 15.8 / 22.9 | 3.89 / 7.91 | 9.25 / 21.8 | 32.3 | 36.4 |

The same build's `scenarios --full` run passed A1–C1 at full size (B6: 105
kill points on a 1,000,000-key database, 237.6 s). It was then stopped by us
because it was an outdated build. The 18 "failures" that log shows from C2
onwards all have exit code 0xC0000142 (process failed to initialise),
starting at the moment the task was stopped. They are artifacts of stopping
it, not results: [scenarios-full-STOPPED.log](results/final/torture/earlier-tester-build-2130/scenarios-full-STOPPED.log).

## 6. Engine evidence from the other suites (same engine code)

| Suite | When | Result |
|---|---|---|
| Go scenario suite, full mode (`KVDB_SCENARIOS=full go test ./src/scenarios`) | 22:18–22:34 | **32 / 32 PASS** (918 s) — [SCENARIO_RESULTS_gotest.md](results/final/SCENARIO_RESULTS_gotest.md) |
| First-generation torture tool, 8 runs (incl. 500 iterations seed 12345) | 19:29–19:45 | all PASS — [results/validation/](results/validation/README.md) |
| 1,000,000 PUT + 1,000,000 DELETE acceptance | 19:45–19:58 | PASS — [results/validation/](results/validation/README.md) |

## 7. What these results do not show

* `torn-tails=0` in almost every run: a real `TerminateProcess` almost never
  lands between the two writes of one frame on this machine. Torn frames are
  shown by the scenario replay of every byte prefix (A2, A7, B1, B2, B6, F4)
  and by the fault-injection suite.
* No power loss and no lying disk: a process kill leaves the OS cache intact.
* One machine, one OS.
* The 11 non-default modes were not re-run with the final tester build. The
  tester changes to `run` since 21:30 are the verification counters, the
  `--keep-going` baseline, the minimizer and failure classification, and not
  sending requests generated after a graceful close (close-reopen mode).
  None of them changes the workload sent to the engine before a kill; the
  results are reported as evidence from the earlier build, as labelled above.
